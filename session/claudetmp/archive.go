package claudetmp

import (
	"archive/zip"
	"compress/flate"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// trashPrefix starts the name of the tombstone Archive renames its source
// to before deleting it.
const trashPrefix = ".loom-trash-"

// ManifestName is the archive entry describing where its content came from.
const ManifestName = ".loom-archive.json"

// Manifest is an archive's ManifestName entry. Restore recreates the
// directory under DirName exactly, which covers a truncated name whose
// hash loom cannot compute.
type Manifest struct {
	DirName       string         `json:"dir_name"`
	Source        string         `json:"source"`
	Worktree      string         `json:"worktree"`
	Reason        string         `json:"reason"`
	ArchivedAt    time.Time      `json:"archived_at"`
	SkippedCaches []SkippedCache `json:"skipped_caches,omitempty"`
}

// SkippedCache is a cache directory Archive left out (slash path relative
// to the archived dir) and the bytes it held.
type SkippedCache struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Result is what one Archive did, for the log.
type Result struct {
	BytesIn    int64 // regular-file bytes archived
	BytesOut   int64 // the zip's size
	CacheBytes int64 // bytes in the cache dirs left out
}

// cacheDirSignature opens every valid CACHEDIR.TAG
// (https://bford.info/cachedir/): cargo's target/, among others.
const cacheDirSignature = "Signature: 8a477f597d28d172789f06886806bc55"

// ArchiveDir is where configDir's archives live.
func ArchiveDir(configDir string) string {
	return filepath.Join(configDir, "archive", "claude-tmp")
}

// archiveFile is what Archive writes the zip through.
type archiveFile interface {
	io.Writer
	Sync() error
	Close() error
}

// openArchiveFile creates the new, empty file (mode 0600) Archive writes,
// <dir>/<pattern> with its last "*" replaced by a random string (see
// os.CreateTemp), and returns it with its name. A var so a test can make
// writing fail half-way.
var openArchiveFile = func(dir, pattern string) (archiveFile, string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	return f, f.Name(), nil
}

// afterZipInPlace runs once Archive has renamed its zip into place and
// before it moves src aside: the window in which another process can act on
// the same src or zip. A var so a test can play that process.
var afterZipInPlace = func() {}

// Archive zips src into zipPath (mode 0600, its dir 0700) and then deletes
// src.
//
// Symlinks are stored as links and never followed (following one could
// archive $HOME). Sockets, FIFOs and devices are skipped, and so is every
// directory holding a valid CACHEDIR.TAG; m.SkippedCaches lists those.
// m.DirName, m.Source and m.ArchivedAt are filled in when empty.
//
// The zip is written to a file of its own beside zipPath,
// "<zip name>.<random>.partial", and renamed into place only once complete
// and synced. The name is never shared, so two processes archiving into one
// directory cannot interleave writes into one file, rename the corrupt
// result into place and delete their sources.
//
// Only after that rename does src go, in two steps: src is renamed to a
// ".loom-trash-<pid>-<nanoseconds>" tombstone in its own directory (atomic,
// so src's name is either whole or gone), and the tombstone is deleted. So
// success means the zip is in place and src's name is gone, and an error
// before the move aside means src is intact and no new zip exists.
//
// If the move aside fails, the zip is removed again, so that src stays whole
// and no new zip is left. The exceptions are where another process may be
// archiving the same src (see abandonZip): a src that is gone, or a zip that
// is no longer the one this call wrote, keeps the zip, and the error says
// so. If removing the zip fails, the error says it is left at its path.
//
// A tombstone that cannot be fully deleted (root-owned files an agent's
// container left, say) is logged and kept: nothing locates or sweeps a
// ".loom-trash-" name.
func Archive(src, zipPath string, m Manifest) (Result, error) {
	if m.DirName == "" {
		m.DirName = filepath.Base(src)
	}
	if m.Source == "" {
		m.Source = src
	}
	if m.ArchivedAt.IsZero() {
		m.ArchivedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create the archive dir: %w", err)
	}
	f, partial, err := openArchiveFile(filepath.Dir(zipPath), filepath.Base(zipPath)+".*.partial")
	if err != nil {
		return Result{}, fmt.Errorf("create a partial file for %s: %w", zipPath, err)
	}
	fail := func(err error) (Result, error) {
		_ = f.Close()
		_ = os.Remove(partial)
		return Result{}, err
	}
	var res Result
	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	if err := addTree(zw, src, &m, &res); err != nil {
		return fail(fmt.Errorf("archive %s: %w", src, err))
	}
	if err := addManifest(zw, m); err != nil {
		return fail(fmt.Errorf("write the manifest: %w", err))
	}
	if err := zw.Close(); err != nil {
		return fail(fmt.Errorf("finish %s: %w", partial, err))
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("sync %s: %w", partial, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partial)
		return Result{}, fmt.Errorf("close %s: %w", partial, err)
	}
	if err := os.Rename(partial, zipPath); err != nil {
		_ = os.Remove(partial)
		return Result{}, fmt.Errorf("move %s into place: %w", zipPath, err)
	}
	written, err := os.Lstat(zipPath) // identifies this call's zip, should it have to be taken back
	if err != nil {
		written = nil
	} else {
		res.BytesOut = written.Size()
	}
	syncDir(filepath.Dir(zipPath))
	afterZipInPlace()
	trash := filepath.Join(filepath.Dir(src), fmt.Sprintf("%s%d-%x", trashPrefix, os.Getpid(), time.Now().UnixNano()))
	if err := os.Rename(src, trash); err != nil {
		return Result{}, abandonZip(zipPath, written, src, err)
	}
	if err := removeTree(trash); err != nil {
		log.For("claudetmp").Warn("claudetmp.trash_kept", "trash", trash, "source", src, "err", err.Error())
	}
	return res, nil
}

// abandonZip decides the fate of the zip Archive just put in place when src
// could not be moved aside (renameErr), and returns the error Archive
// reports. Another process archiving the same src can be at work, so the
// zip is removed only when it is certainly this call's and src certainly
// still exists:
//
//   - src is gone (renameErr is ErrNotExist): someone else moved or deleted
//     it, so the zip is a complete copy, perhaps the only one. Kept.
//   - the file at zipPath is not the one this call wrote (written, from just
//     after its own rename): another process's zip replaced it. Kept. So is
//     a zip whose identity could not be checked.
//   - otherwise the zip is removed again, so that an error leaves src whole
//     and no new zip behind. If that fails, the error says the zip is left.
//
// The check and the removal are two steps, so a replacement between them
// can still be lost; the window is a few microseconds, not an archive.
func abandonZip(zipPath string, written fs.FileInfo, src string, renameErr error) error {
	if errors.Is(renameErr, fs.ErrNotExist) {
		return fmt.Errorf("move %s aside: %w (it is gone, so another process moved it; the archive %s is kept)", src, renameErr, zipPath)
	}
	cur, err := os.Lstat(zipPath)
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("move %s aside: %w", src, renameErr) // already gone: nothing to take back
	case err != nil || written == nil || !os.SameFile(written, cur):
		return fmt.Errorf("move %s aside: %w (the archive %s is kept: it is not certainly the one this call wrote)", src, renameErr, zipPath)
	}
	if rmErr := os.Remove(zipPath); rmErr != nil {
		return fmt.Errorf("move %s aside: %w (the archive %s was written and could not be removed: %v)", src, renameErr, zipPath, rmErr)
	}
	return fmt.Errorf("move %s aside: %w", src, renameErr)
}

// syncDir fsyncs dir so a rename into it survives a crash. Best effort:
// the zip is complete either way, and a failure here changes nothing a
// caller could act on.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// addTree adds src's content to zw, named by slash paths relative to src.
// WalkDir never follows symlinks.
func addTree(zw *zip.Writer, src string, m *Manifest, res *Result) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime()}
			hdr.SetMode(mode)
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, target)
			return err
		case mode.IsDir():
			if isCacheDir(p) {
				n := treeSize(p)
				m.SkippedCaches = append(m.SkippedCaches, SkippedCache{Path: name, Bytes: n})
				res.CacheBytes += n
				return fs.SkipDir
			}
			hdr := &zip.FileHeader{Name: name + "/", Modified: info.ModTime()}
			hdr.SetMode(mode)
			_, err := zw.CreateHeader(hdr)
			return err
		case mode.IsRegular():
			return addFile(zw, p, name, info, res)
		default: // sockets, FIFOs, devices
			return nil
		}
	})
}

// addFile adds the regular file p. It checks the opened file is still the
// one WalkDir saw, so a file swapped for a symlink since is never followed.
func addFile(zw *zip.Writer, p, name string, info fs.FileInfo, res *Result) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !os.SameFile(info, fi) {
		return fmt.Errorf("%s changed while being archived", p)
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, f)
	res.BytesIn += n
	return err
}

// addManifest writes m as the archive's ManifestName entry.
func addManifest(zw *zip.Writer, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: m.ArchivedAt}
	hdr.SetMode(0o600)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// isCacheDir reports whether dir holds a regular CACHEDIR.TAG that starts
// with the spec's signature. A malformed tag is ordinary content.
func isCacheDir(dir string) bool {
	tag := filepath.Join(dir, "CACHEDIR.TAG")
	if fi, err := os.Lstat(tag); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(tag)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(cacheDirSignature))
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == cacheDirSignature
}

// treeSize sums the regular files under dir, for the log.
func treeSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// removeTree deletes dir. A tree with read-only directories (Go's module
// cache makes its own so) refuses RemoveAll, so on failure it gives the
// owner full permission on every directory and tries once more. WalkDir
// visits a directory before reading it, so the chmod lands in time.
func removeTree(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil {
				_ = os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// PurgeTrash removes what an interrupted Archive or Restore left under root:
// every directory directly under root whose name starts with ".loom-trash-"
// (an Archive tombstone) or ".loom-restore-" (a Restore's staging directory)
// and whose own modification time is more than olderThan ago. It returns how
// many it removed. A missing or unreadable root removes nothing.
//
// Neither holds anything that is not safe elsewhere. Archive renames its
// source to a tombstone only after the zip is in place, so a tombstone holds
// content already archived; one survives when a quit or takeover interrupted
// the delete, or the delete failed. Restore extracts into a staging
// directory and deletes its zip only after renaming that into place, so a
// staging directory left by a quit, takeover or crash is a partial copy of
// a zip that still exists. Nothing else would retry either.
//
// Removal is best effort; failures are logged at debug. The time is the
// directory's own mtime, which moves only when its direct entries do, and
// which the rename into a tombstone leaves as it was. So a tombstone can
// look old the moment it is made, which is harmless: racing the delete that
// follows only removes what is already doomed. A staging directory can look
// old while a restore is still filling its subtree, so olderThan must
// exceed the longest plausible restore (the sweep uses an hour). Past that,
// a purge racing the restore's rename can keep deleting inside what has
// just become the restored dir (RemoveAll works through the fd it opened),
// and Restore then deletes the zip. Only directories with these
// prefixes are touched: never a session's temp dir, however old.
func PurgeTrash(root string, olderThan time.Duration) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !(strings.HasPrefix(e.Name(), trashPrefix) || strings.HasPrefix(e.Name(), stagingPrefix)) {
			continue
		}
		p := filepath.Join(root, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || !fi.ModTime().Before(cutoff) {
			continue
		}
		if err := removeTree(p); err != nil {
			log.For("claudetmp").Debug("claudetmp.trash_purge_failed", "trash", p, "err", err.Error())
			continue
		}
		removed++
	}
	return removed
}

// PurgePartials removes the partial files Archive left in dir: every regular
// file directly in dir whose name ends in ".partial" and whose modification
// time is more than olderThan ago. It returns how many it removed. A
// missing or unreadable dir removes nothing.
//
// Each Archive writes to a uniquely named partial file (see Archive), so one
// a crash or a quit left behind is never overwritten by a retry, and
// nothing else removes it. It holds an incomplete zip and nothing that is
// not still in its source, which is only deleted after the zip is complete.
// A live archive keeps writing, which keeps its mtime fresh, so olderThan
// need only be longer than any pause in writing; the sweep uses 24 hours.
// Removal is best effort; failures are logged at debug. Directories and
// files without the suffix are never touched, zips included.
func PurgePartials(dir string, olderThan time.Duration) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".partial") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(p); err != nil {
			log.For("claudetmp").Debug("claudetmp.partial_purge_failed", "partial", p, "err", err.Error())
			continue
		}
		removed++
	}
	return removed
}

// ArchiveAs archives src (see Archive) as <dir>/<name>.zip and returns
// that path. An existing <name>.zip, parked by a pause whose restore later
// failed, is first renamed to <name>.<UTC timestamp>.zip: a permanent
// archive that no resume restores. The timestamp is the earlier zip's own
// modification time, about when it was archived, rather than when it was
// moved aside.
func ArchiveAs(dir, name, src string, m Manifest) (string, Result, error) {
	zipPath := filepath.Join(dir, name+".zip")
	if fi, err := os.Lstat(zipPath); err == nil {
		stamp := fi.ModTime()
		if stamp.IsZero() {
			stamp = time.Now()
		}
		if err := os.Rename(zipPath, demotedPath(dir, name, stamp)); err != nil {
			return zipPath, Result{}, fmt.Errorf("move the earlier %s aside: %w", zipPath, err)
		}
	}
	res, err := Archive(src, zipPath, m)
	return zipPath, res, err
}

// demotedPath is a free <dir>/<name>.<UTC timestamp of at>[-N].zip.
func demotedPath(dir, name string, at time.Time) string {
	stamp := at.UTC().Format("20060102T150405Z")
	p := filepath.Join(dir, fmt.Sprintf("%s.%s.zip", name, stamp))
	for n := 2; ; n++ {
		if _, err := os.Lstat(p); err != nil {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s.%s-%d.zip", name, stamp, n))
	}
}

// Parked returns the zip a pause parked for the session whose cwd was
// worktreePath: <dir>/<name>.zip, where name is ArchiveName of one of
// Names(worktreePath). Demoted archives carry a "." in their name and
// never match, and neither does a truncated name (see Name.Matches): such a
// session's temp dir is never archived, so no zip of it is parked.
func Parked(dir, worktreePath string, prefixes []string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var wants []Name
	for _, n := range Names(worktreePath) {
		wants = append(wants, Name{Value: ArchiveName(n.Value, prefixes), Truncated: n.Truncated})
	}
	var found []string
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ".zip")
		if !ok || strings.Contains(base, ".") || !e.Type().IsRegular() {
			continue
		}
		for _, w := range wants {
			if w.Matches(base) {
				found = append(found, e.Name())
				break
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return filepath.Join(dir, found[0]), true
}

// DirSize sums the sizes of the regular files directly in dir: the archive
// dir's total, for the log.
func DirSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}
