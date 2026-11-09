package flowview

import (
	"path/filepath"
	"regexp"
)

var notKey = regexp.MustCompile(`[^A-Za-z0-9]`)

// RepoKey is the repo key flowconf.repo_key prints (`loom flow conf key`):
// the main checkout's resolved path (the parent of git's common dir, or
// the common dir itself for a bare repository) with every character
// outside [A-Za-z0-9] made -. Records live under <records>/<key>/.
func RepoKey(worktree string) (string, error) {
	common, err := gitOut(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	main := common
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	if resolved, err := filepath.EvalSymlinks(main); err == nil {
		main = resolved
	}
	return notKey.ReplaceAllString(main, "-"), nil
}
