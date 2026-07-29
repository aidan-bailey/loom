package review

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const (
	critDir    = ".crit"
	reviewsDir = "reviews"
)

// EnsureDirs creates <root>/.crit/reviews and a self-ignoring
// .gitignore so review state never shows up in git status or loom's
// diff stats.
func EnsureDirs(root string) error {
	dir := filepath.Join(root, critDir, reviewsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	gitignorePath := filepath.Join(root, critDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("creating .crit/.gitignore: %w", err)
		}
	}
	return nil
}

// ReviewPath maps a document path to its review file under root. The
// hash covers the absolute doc path so renaming the worktree parent
// (loom never does) is the only way to orphan a review.
func ReviewPath(root, docPath string) string {
	abs, err := filepath.Abs(docPath)
	if err != nil {
		abs = docPath
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(abs)))
	return filepath.Join(root, critDir, reviewsDir, hash+".yaml")
}
