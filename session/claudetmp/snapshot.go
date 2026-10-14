package claudetmp

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// ChatArchiveDir is where configDir's chat archives live by default.
func ChatArchiveDir(configDir string) string {
	return filepath.Join(configDir, "archive", "chats")
}

// snapshotSlack is how long before its walk began a snapshot's zip is
// stamped. File times come from a coarse clock (a kernel tick, or whole
// seconds on some filesystems), so a write during the walk can carry a time
// slightly before the walk's start; stamping the zip earlier still makes
// every such write read as newer at the next Snapshot. The cost is at most
// one redundant snapshot of a file written just before a walk.
const snapshotSlack = 2 * time.Second

// Snapshot copies src, a Claude project dir holding a session's chats, into
// zipPath (mode 0600, its dir 0700), and never touches src. It reports
// whether it wrote a zip.
//
// When zipPath exists and nothing under src (src included) was modified
// after the zip's own mtime, it writes nothing. Otherwise it writes a fresh
// zip: src's content, walked as Archive walks it, then every entry of the
// previous zip that src no longer has (Claude's cleanup deleted the file),
// copied across without recompression and named in m.CarriedForward. A
// file on disk always replaces its archived copy, and nothing an earlier
// snapshot held is ever dropped. m.DirName, m.Source and m.ArchivedAt are
// filled in when empty.
//
// The zip is written to a partial file of its own and renamed into place
// once complete and synced, as Archive does; its mtime is then set to
// snapshotSlack before the walk began. A previous zip that cannot be read
// is moved aside to <name>.<UTC timestamp>.zip and kept, and the new zip
// starts fresh. On any error the partial file is removed and the previous
// zip is left as it was (or where it was moved).
func Snapshot(src, zipPath string, m Manifest) (Result, bool, error) {
	start := time.Now()
	if fi, err := os.Lstat(src); err != nil {
		return Result{}, false, fmt.Errorf("read %s: %w", src, err)
	} else if !fi.IsDir() {
		return Result{}, false, fmt.Errorf("%s is not a directory", src)
	}
	dir := filepath.Dir(zipPath)
	var prev *zip.ReadCloser
	if zi, err := os.Lstat(zipPath); err == nil {
		if !modifiedSince(src, zi.ModTime()) {
			return Result{}, false, nil
		}
		if prev, err = zip.OpenReader(zipPath); err != nil {
			prev = nil
			name := strings.TrimSuffix(filepath.Base(zipPath), ".zip")
			if err := os.Rename(zipPath, demotedPath(dir, name, zi.ModTime())); err != nil {
				return Result{}, false, fmt.Errorf("move the unreadable %s aside: %w", zipPath, err)
			}
		}
	}
	closePrev := func() {
		if prev != nil {
			_ = prev.Close()
			prev = nil
		}
	}
	defer closePrev()

	if m.DirName == "" {
		m.DirName = filepath.Base(src)
	}
	if m.Source == "" {
		m.Source = src
	}
	if m.ArchivedAt.IsZero() {
		m.ArchivedAt = start.UTC()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, false, fmt.Errorf("create the archive dir: %w", err)
	}
	f, partial, err := openArchiveFile(dir, filepath.Base(zipPath)+".*.partial")
	if err != nil {
		return Result{}, false, fmt.Errorf("create a partial file for %s: %w", zipPath, err)
	}
	fail := func(err error) (Result, bool, error) {
		_ = f.Close()
		_ = os.Remove(partial)
		return Result{}, false, err
	}
	var res Result
	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	seen := map[string]bool{}
	if err := addTree(zw, src, &m, &res, seen); err != nil {
		return fail(fmt.Errorf("archive %s: %w", src, err))
	}
	if prev != nil {
		for _, e := range prev.File {
			if e.Name == ManifestName || seen[e.Name] {
				continue
			}
			if err := zw.Copy(e); err != nil {
				return fail(fmt.Errorf("carry %q forward: %w", e.Name, err))
			}
			seen[e.Name] = true
			m.CarriedForward = append(m.CarriedForward, e.Name)
		}
	}
	res.Carried = len(m.CarriedForward)
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
		return Result{}, false, fmt.Errorf("close %s: %w", partial, err)
	}
	closePrev() // Windows renames over no open file
	if err := os.Rename(partial, zipPath); err != nil {
		_ = os.Remove(partial)
		return Result{}, false, fmt.Errorf("move %s into place: %w", zipPath, err)
	}
	syncDir(dir)
	stamp := start.Add(-snapshotSlack)
	if err := os.Chtimes(zipPath, stamp, stamp); err != nil {
		// The zip is complete. Its rename time stands in, so a write during
		// the walk may wait for the next change to be copied.
		log.For("claudetmp").Warn("claudetmp.snapshot_stamp_failed", "zip", zipPath, "err", err.Error())
	}
	if fi, err := os.Lstat(zipPath); err == nil {
		res.BytesOut = fi.Size()
	}
	return res, true, nil
}

// modifiedSince reports whether dir or anything under it was modified after
// t. A deletion counts: it changes its directory's mtime. It fails open: an
// entry it cannot read counts as modified, so the snapshot that follows
// reports the error.
func modifiedSince(dir string, t time.Time) bool {
	changed := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(t) {
			changed = true
			return fs.SkipAll
		}
		return nil
	})
	return changed || err != nil
}

// Fill extracts into dir every entry of the archive at zipPath that dir
// lacks, and returns how many files and links it wrote. It never
// overwrites, never deletes the zip, and leaves what it writes with the
// current time as its mtime, so Claude's age-based cleanup does not take a
// restored transcript straight away. dir's base name must be the
// manifest's dir_name: the archive holds that dir's content and no other's.
// dir is created (0700) when missing.
//
// Entries are written through an os.Root, which rejects "..", absolute
// paths and escapes through symlinks. An error stops it part way, and what
// it already wrote stays: it only filled gaps.
//
// No error names zipPath: the caller does (see Restore).
func Fill(zipPath, dir string) (int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return 0, fmt.Errorf("open the archive: %w", err)
	}
	defer zr.Close()
	manifest, err := manifestEntry(&zr.Reader)
	if err != nil {
		return 0, err
	}
	m, err := readManifest(manifest)
	if err != nil {
		return 0, err
	}
	if m.DirName != filepath.Base(dir) {
		return 0, fmt.Errorf("the archive holds %q, not %q", m.DirName, filepath.Base(dir))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("create %s: %w", dir, err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	n := 0
	for _, f := range zr.File {
		if f == manifest {
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || name == "." {
			return n, fmt.Errorf("archive entry %q is not a relative path inside the archive", f.Name)
		}
		if _, err := r.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return n, fmt.Errorf("restore %q: %w", f.Name, err)
		}
		if parent := path.Dir(name); parent != "." {
			if err := r.MkdirAll(parent, 0o700); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
		}
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := readEntry(f, 4096)
			if err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			if err := r.Symlink(string(target), name); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			n++
		case mode.IsDir():
			if err := r.MkdirAll(name, 0o700); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
		case mode.IsRegular():
			if err := writeEntry(r, f, name); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			if err := r.Chmod(name, mode.Perm()); err != nil {
				return n, fmt.Errorf("restore the mode of %q: %w", f.Name, err)
			}
			n++
		}
	}
	return n, nil
}
