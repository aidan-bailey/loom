package flowview

import (
	"slices"
	"strings"
)

// branchFiles is every path the branch's own commits since base touched
// (git log --first-parent --no-merges --name-only base..HEAD: a merge of
// main brings in none of main's files) and every path git status reports,
// untracked files included, relative to top, sorted, without duplicates.
// A base git doesn't know is an error.
func branchFiles(top, base string) ([]string, error) {
	logged, err := gitRaw(top, "log", "-z", "--first-parent", "--no-merges", "--name-only", "--format=", base+"..HEAD", "--")
	if err != nil {
		return nil, err
	}
	// --no-optional-locks: a status refreshes the index otherwise, and a
	// reader writes nothing.
	status, err := gitRaw(top, "--no-optional-locks", "status", "-z", "--porcelain", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(logged, "\x00") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	for _, entry := range strings.Split(status, "\x00") {
		if len(entry) > 3 {
			files = append(files, entry[3:])
		}
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// headTree is which of paths HEAD's tree holds (git ls-tree), relative to
// top. A repository with no commit yet holds none.
func headTree(top string, paths []string) (map[string]bool, error) {
	in := map[string]bool{}
	if len(paths) == 0 {
		return in, nil
	}
	if _, err := gitOut(top, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return in, nil
	}
	out, err := gitRaw(top, append([]string{"ls-tree", "-z", "-r", "--name-only", "HEAD", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			in[f] = true
		}
	}
	return in, nil
}
