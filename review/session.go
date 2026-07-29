package review

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CodeReviewSession tracks which files belong to the current code
// review. Written for interop: an agent running the crit CLI in the
// worktree reads the same manifest.
type CodeReviewSession struct {
	Files     []string  `yaml:"files"`
	DiffBase  string    `yaml:"diff_base"`
	CreatedAt time.Time `yaml:"created_at"`
}

func sessionPath(root string) string {
	return filepath.Join(root, critDir, "code-review.yaml")
}

func SaveSession(root string, session *CodeReviewSession) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	data, err := yaml.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	if err := os.WriteFile(sessionPath(root), data, 0o644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}

func LoadSession(root string) (*CodeReviewSession, error) {
	data, err := os.ReadFile(sessionPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no active code review session")
		}
		return nil, fmt.Errorf("reading session: %w", err)
	}
	var session CodeReviewSession
	if err := yaml.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing session: %w", err)
	}
	return &session, nil
}
