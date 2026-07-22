package files

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// mdSkipDirs are directory names pruned from the markdown follow scan.
// Broader than listViaWalk's pruning because this walk runs every
// health tick: dependency and build trees are both noisy (their .md
// files are never the user's working documents) and large.
var mdSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".loom":        true,
	"dist":         true,
	"target":       true,
}

// MostRecentMarkdown walks root and returns the most recently modified
// markdown file (.md/.markdown, case-insensitive). ok=false when the
// tree holds none. Unreadable entries and symlinks are skipped rather
// than failing the scan — a permission error on one subtree must not
// blank the follow view. Callers run this inside a tea.Cmd, so a slow
// filesystem stalls one background goroutine, not the UI loop.
func MostRecentMarkdown(root string) (path string, mtime time.Time, ok bool, err error) {
	if root == "" {
		return "", time.Time{}, false, errors.New("files.MostRecentMarkdown: root is empty")
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && mdSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if !ok || info.ModTime().After(mtime) {
			path, mtime, ok = p, info.ModTime(), true
		}
		return nil
	})
	if err != nil {
		return "", time.Time{}, false, err
	}
	return path, mtime, ok, nil
}
