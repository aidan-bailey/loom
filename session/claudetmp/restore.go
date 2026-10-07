package claudetmp

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aidan-bailey/loom/log"
)

// stagingPrefix starts the name of the sibling directory Restore extracts
// into before renaming it into place.
const stagingPrefix = ".loom-restore-"

// Restore recreates the temp dir an archive holds under root, named by the
// manifest's exact dir_name, then deletes the zip. root is created (0700)
// when missing: /tmp is often tmpfs, which a reboot empties.
//
// It refuses when that directory already exists. Entries are extracted into
// a fresh sibling through an os.Root, which rejects "..", absolute paths and
// escapes through symlinks, and the sibling is renamed into place only once
// complete. On any error the sibling is removed and the zip kept.
//
// No error names zipPath: the caller does, and an error bar truncates a
// message that repeats a long path. (os.Open's own error carries the path,
// so a failed open is reported by its cause alone.)
func Restore(zipPath, root string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("open the archive: %w", err)
	}
	err = restore(&zr.Reader, root)
	_ = zr.Close()
	if err != nil {
		return err
	}
	if err := os.Remove(zipPath); err != nil {
		log.For("claudetmp").Warn("claudetmp.restored_zip_kept", "zip", zipPath, "err", err.Error())
	}
	return nil
}

// restore is Restore with the zip open.
func restore(zr *zip.Reader, root string) error {
	manifest, err := manifestEntry(zr)
	if err != nil {
		return err
	}
	m, err := readManifest(manifest)
	if err != nil {
		return err
	}
	if !fs.ValidPath(m.DirName) || m.DirName == "." || strings.ContainsAny(m.DirName, `/\`) {
		return fmt.Errorf("the archive's manifest names an invalid directory %q", m.DirName)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	target := filepath.Join(root, m.DirName)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%q already exists", target)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %q: %w", target, err)
	}
	staging, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		return fmt.Errorf("create a staging dir under %s: %w", root, err)
	}
	if err := extract(zr, staging, manifest); err != nil {
		_ = removeTree(staging)
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = removeTree(staging)
		return fmt.Errorf("move the restored dir into place at %q: %w", target, err)
	}
	return nil
}

// manifestEntry returns the archive's ManifestName entry: the last one of
// that name, which Archive always writes last, so a user file of the same
// name at the top level of the archived dir cannot stand in for it.
func manifestEntry(zr *zip.Reader) (*zip.File, error) {
	for i := len(zr.File) - 1; i >= 0; i-- {
		if zr.File[i].Name == ManifestName {
			return zr.File[i], nil
		}
	}
	return nil, fmt.Errorf("the archive has no %s", ManifestName)
}

// readManifest decodes the manifest entry f.
func readManifest(f *zip.File) (Manifest, error) {
	var m Manifest
	rc, err := f.Open()
	if err != nil {
		return m, fmt.Errorf("read the archive's manifest: %w", err)
	}
	defer rc.Close()
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("parse the archive's manifest: %w", err)
	}
	return m, nil
}

// extract writes zr's entries, all but the manifest entry, under dir through
// an os.Root, so no entry can land outside it. Modes are applied last and
// deepest first, so a read-only directory does not block its own content.
func extract(zr *zip.Reader, dir string, manifest *zip.File) error {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	type pendingMode struct {
		name string
		perm fs.FileMode
	}
	var modes []pendingMode
	for _, f := range zr.File {
		if f == manifest {
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || name == "." {
			return fmt.Errorf("archive entry %q is not a relative path inside the archive", f.Name)
		}
		if parent := path.Dir(name); parent != "." {
			if err := r.MkdirAll(parent, 0o700); err != nil {
				return fmt.Errorf("restore %q: %w", f.Name, err)
			}
		}
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := readEntry(f, 4096)
			if err != nil {
				return fmt.Errorf("restore %q: %w", f.Name, err)
			}
			if err := r.Symlink(string(target), name); err != nil {
				return fmt.Errorf("restore %q: %w", f.Name, err)
			}
		case mode.IsDir():
			if err := r.MkdirAll(name, 0o700); err != nil {
				return fmt.Errorf("restore %q: %w", f.Name, err)
			}
			modes = append(modes, pendingMode{name, mode.Perm()})
		case mode.IsRegular():
			if err := writeEntry(r, f, name); err != nil {
				return fmt.Errorf("restore %q: %w", f.Name, err)
			}
			modes = append(modes, pendingMode{name, mode.Perm()})
		}
	}
	for i := len(modes) - 1; i >= 0; i-- {
		if err := r.Chmod(modes[i].name, modes[i].perm); err != nil {
			return fmt.Errorf("restore the mode of %q: %w", modes[i].name, err)
		}
	}
	return nil
}

// writeEntry writes the regular-file entry f as name inside r.
func writeEntry(r *os.Root, f *zip.File, name string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// readEntry reads at most limit bytes of entry f: a symlink's target.
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}
