package review

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// Load reads the review state for docPath, returning an empty state
// when none exists yet.
func Load(root, docPath string) (*ReviewState, error) {
	data, err := os.ReadFile(ReviewPath(root, docPath))
	if err != nil {
		if os.IsNotExist(err) {
			return &ReviewState{File: docPath, Comments: []Comment{}}, nil
		}
		return nil, fmt.Errorf("reading review: %w", err)
	}
	var state ReviewState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing review YAML: %w", err)
	}
	return &state, nil
}

// Save writes state atomically (tmp + rename) under a flock so a
// concurrently running crit CLI in the same worktree can't interleave.
func Save(root string, state *ReviewState) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	reviewPath := ReviewPath(root, state.File)
	lockPath := reviewPath + ".lock"

	fileLock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring review lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("review file %s is locked by another process", lockPath)
	}
	defer func() {
		_ = fileLock.Unlock()
		_ = os.Remove(lockPath)
	}()

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling review: %w", err)
	}
	tmpPath := reviewPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing temp review file: %w", err)
	}
	if err := os.Rename(tmpPath, reviewPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp review file: %w", err)
	}
	return nil
}
