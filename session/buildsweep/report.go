// Package buildsweep deletes the Rust executables in session worktrees'
// target dirs that have gone unused for a while: cargo writes a new test
// binary under a new hash for each feature set and profile, and never
// deletes the old one. It reads only the filesystem, /proc and the mount
// table; the model (core) decides which sessions to give it.
package buildsweep

import "time"

// Options are a sweep request's settings. DryRun reports what would go
// and deletes nothing; AfterHours, when not 0, replaces the
// build_sweep_after_hours setting for this sweep.
type Options struct {
	DryRun     bool
	AfterHours int
}

// Skip names why a session's worktree was not swept; "" when it was.
type Skip string

// The reasons a worktree is skipped, as the report and the log name them.
const (
	// SkipStatus: the session's agent is not idle (the model's call).
	SkipStatus Skip = "status"
	// SkipNoTarget: the worktree has no cargo target dir.
	SkipNoTarget Skip = "no_target"
	// SkipAtimeOff: its filesystem records no access times, or too
	// coarsely for the threshold, or they can't be checked.
	SkipAtimeOff Skip = "atime_off"
	// SkipBuilding: cargo holds one of the target dir's locks.
	SkipBuilding Skip = "building"
	// SkipTesting: a cargo process runs in the worktree.
	SkipTesting Skip = "testing"
	// SkipError: the worktree or its target dir could not be read.
	SkipError Skip = "error"
)

// Target is one session's worktree, named as the report shows it. One
// whose Skip is set is reported as given and its disk never read.
type Target struct {
	Workspace string
	Title     string
	Status    string
	Worktree  string
	Skip      Skip
	Detail    string
}

// File is one executable the sweep deleted, or would in a dry run, with
// the time it was last used (its access or modification time, the later).
type File struct {
	Path     string // relative to the worktree
	Size     int64
	LastUsed time.Time
}

// Result is one session's sweep: its Target, with Skip and Detail set
// when it was skipped, and otherwise the files that went, the bytes they
// held and the deletes that failed.
type Result struct {
	Target
	Files  []File
	Freed  int64
	Errors []string
}

// Report is one sweep over every target it was given.
type Report struct {
	DryRun     bool
	AfterHours int
	Results    []Result
}
