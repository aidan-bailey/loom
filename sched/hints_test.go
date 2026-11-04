package sched

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHints_AddsWhatTheEnvironmentLacks(t *testing.T) {
	got := Hints([]string{"PATH=/bin", "HOME=/home/me"}, 4, 14)
	assert.Equal(t, []string{
		"PATH=/bin",
		"HOME=/home/me",
		"CARGO_BUILD_JOBS=4",
		"RUST_TEST_THREADS=4",
		"MAKEFLAGS=-j4",
		"NIX_BUILD_CORES=4",
		"CMAKE_BUILD_PARALLEL_LEVEL=4",
		"GOFLAGS=-p=4",
		"LOOM_JOB=14",
	}, got)

	assert.Len(t, Hints(nil, 2, 1), 7, "an empty environment gets the six hints and LOOM_JOB")
}

// TestHints_KeepsWhatTheSubmitterSet: a variable the submitter's environment
// already sets keeps its value, an empty one included, and is not repeated; a
// name that only starts like a hint is no hint.
func TestHints_KeepsWhatTheSubmitterSet(t *testing.T) {
	env := []string{"CARGO_BUILD_JOBS=2", "GOFLAGS=-mod=vendor", "MAKEFLAGS=", "CARGO_BUILD_JOBS_EXTRA=1"}
	got := Hints(env, 8, 3)
	assert.Equal(t, []string{
		"CARGO_BUILD_JOBS=2",
		"GOFLAGS=-mod=vendor",
		"MAKEFLAGS=",
		"CARGO_BUILD_JOBS_EXTRA=1",
		"RUST_TEST_THREADS=8",
		"NIX_BUILD_CORES=8",
		"CMAKE_BUILD_PARALLEL_LEVEL=8",
		"LOOM_JOB=3",
	}, got)
}

// TestHints_ReplacesAnInheritedLoomJob: a job submitted from inside a job
// inherits its LOOM_JOB, and the new job's own replaces it, wherever it sat.
func TestHints_ReplacesAnInheritedLoomJob(t *testing.T) {
	got := Hints([]string{"A=1", "LOOM_JOB=9", "B=2"}, 2, 10)
	assert.Equal(t, []string{"A=1", "B=2"}, got[:2])
	assert.Equal(t, "LOOM_JOB=10", got[len(got)-1])
	loomJobs := 0
	for _, e := range got {
		if strings.HasPrefix(e, "LOOM_JOB=") {
			loomJobs++
		}
	}
	assert.Equal(t, 1, loomJobs, "exactly one LOOM_JOB")
}

// TestHints_LeavesItsInputAlone: the result is a new slice, and Hints writes
// nothing into the caller's spare capacity.
func TestHints_LeavesItsInputAlone(t *testing.T) {
	env := make([]string, 2, 16)
	env[0], env[1] = "A=1", "LOOM_JOB=9"
	before := slices.Clone(env)
	_ = Hints(env, 2, 3)
	assert.Equal(t, before, env)
	for _, spare := range env[len(env):cap(env)] {
		assert.Empty(t, spare, "Hints wrote into the caller's spare capacity")
	}
}
