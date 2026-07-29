package gitdiff

// Package gitdiff parses git diffs into per-file changed-line maps for
// the review tab. Derived from kevindutra/crit internal/git (see
// NOTICE.md); modified to run against an explicit repo dir via `git -C`
// instead of the process working directory. Kept separate from
// session/git, which shells out for worktree lifecycle operations.

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// ChangeStatus represents the type of change to a file.
type ChangeStatus int

const (
	StatusModified ChangeStatus = iota
	StatusAdded
	StatusDeleted
	StatusRenamed
	StatusUntracked
	StatusBinary
)

// FileChange describes a single changed file.
type FileChange struct {
	Path    string
	Status  ChangeStatus
	OldPath string // for renames
}

// ChangedFiles returns files with changes relative to HEAD (staged + unstaged + untracked).
func ChangedFiles(dir string) ([]FileChange, error) {
	return ChangedFilesFrom(dir, "HEAD")
}

// ChangedFilesFrom returns files changed relative to a ref (branch, commit, HEAD~N).
func ChangedFilesFrom(dir, ref string) ([]FileChange, error) {
	files, err := diffNameStatus(dir, ref)
	if err != nil {
		return nil, err
	}

	// Detect binary files via --numstat (binary shows - - for counts)
	binaries, err := detectBinaryFiles(dir, ref)
	if err != nil {
		return nil, err
	}

	for i := range files {
		if binaries[files[i].Path] {
			files[i].Status = StatusBinary
		}
	}

	// Add untracked files (only for HEAD-based diffs)
	if ref == "HEAD" {
		untracked, err := untrackedFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range untracked {
			files = append(files, FileChange{
				Path:   path,
				Status: StatusUntracked,
			})
		}
	}

	return files, nil
}

// DiffInfo contains parsed diff information for a single file.
type DiffInfo struct {
	ChangedLines map[int]bool          // added/modified line numbers (1-based) in new file
	DeletedAfter map[int][]DeletedLine // deleted lines keyed by the new-file line they appear after (0 = before line 1)
}

// DeletedLine represents a line that was deleted from the old version.
type DeletedLine struct {
	OldLineNum int
	Content    string
}

// DiffFile returns full diff information for a file relative to the given ref.
func DiffFile(dir, path, ref string) (*DiffInfo, error) {
	out, err := gitCommand(dir, "diff", ref, "--", path)
	if err != nil {
		return nil, fmt.Errorf("git diff for %s: %w", path, err)
	}

	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	files, _, err := gitdiff.Parse(strings.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("parsing diff for %s: %w", path, err)
	}

	info := &DiffInfo{
		ChangedLines: make(map[int]bool),
		DeletedAfter: make(map[int][]DeletedLine),
	}

	for _, f := range files {
		for _, frag := range f.TextFragments {
			newLine := frag.NewPosition
			oldLine := frag.OldPosition
			// Track the last new-file line we've seen (for anchoring deletions)
			lastNewLine := int(newLine) - 1 // before the hunk starts

			for _, line := range frag.Lines {
				switch line.Op {
				case gitdiff.OpAdd:
					info.ChangedLines[int(newLine)] = true
					lastNewLine = int(newLine)
					newLine++
				case gitdiff.OpContext:
					lastNewLine = int(newLine)
					newLine++
					oldLine++
				case gitdiff.OpDelete:
					content := strings.TrimSuffix(line.Line, "\n")
					info.DeletedAfter[lastNewLine] = append(info.DeletedAfter[lastNewLine], DeletedLine{
						OldLineNum: int(oldLine),
						Content:    content,
					})
					oldLine++
				}
			}
		}
	}

	return info, nil
}

// diffNameStatus runs git diff --name-status and parses the output.
func diffNameStatus(dir, ref string) ([]FileChange, error) {
	out, err := gitCommand(dir, "diff", ref, "--name-status")
	if err != nil {
		return nil, fmt.Errorf("git diff --name-status: %w", err)
	}

	var files []FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		status := parts[0]
		path := parts[1]

		fc := FileChange{Path: path}
		switch {
		case status == "M":
			fc.Status = StatusModified
		case status == "A":
			fc.Status = StatusAdded
		case status == "D":
			fc.Status = StatusDeleted
		case strings.HasPrefix(status, "R"):
			fc.Status = StatusRenamed
			if len(parts) >= 3 {
				fc.OldPath = parts[1]
				fc.Path = parts[2]
			}
		default:
			fc.Status = StatusModified
		}

		files = append(files, fc)
	}

	return files, nil
}

// detectBinaryFiles returns a set of paths that are binary.
func detectBinaryFiles(dir, ref string) (map[string]bool, error) {
	out, err := gitCommand(dir, "diff", ref, "--numstat")
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat: %w", err)
	}

	binaries := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// Binary files show "-\t-\tpath"
		if strings.HasPrefix(line, "-\t-\t") {
			path := strings.TrimPrefix(line, "-\t-\t")
			binaries[path] = true
		}
	}

	return binaries, nil
}

// untrackedFiles returns untracked file paths.
func untrackedFiles(dir string) ([]string, error) {
	out, err := gitCommand(dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// gitCommand runs a git command and returns stdout.
func gitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// IsGitRepo checks if the given directory is inside a git repository.
func IsGitRepo(dir string) bool {
	_, err := gitCommand(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}
