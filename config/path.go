package config

import "path/filepath"

// CanonicalPath is p with its symlinks resolved, so two spellings of one
// directory compare equal. A path that no longer exists (a deleted
// worktree) resolves through its nearest existing ancestor. Returns "" for
// an empty or relative path.
func CanonicalPath(p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
