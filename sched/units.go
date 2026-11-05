package sched

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// unitTag is the 8 hex digits that keep two looms' units apart under one
// user manager (cgroup spec §15, 1): the SHA-256 of the jobs directory,
// cleaned.
func unitTag(jobsDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(jobsDir)))
	return hex.EncodeToString(sum[:4])
}

// SliceName is the slice the jobs of the loom whose jobs directory is
// jobsDir run in: loom_<tag>.slice, directly under the user's manager beside
// the agents' panes. A dash would nest it in loom.slice, whose weight would
// then be all the panes see of the jobs.
func SliceName(jobsDir string) string { return "loom_" + unitTag(jobsDir) + ".slice" }

// ScopeName is job id's scope for its attempt (0 for its first run):
// loom-<tag>-job-<id>-<attempt+1>.scope.
func ScopeName(jobsDir string, id JobID, attempt int) string {
	return fmt.Sprintf("loom-%s-job-%d-%d.scope", unitTag(jobsDir), id, attempt+1)
}

// CPUWeight is a job's scope weight: 100 per claimed core, 1–10000.
func CPUWeight(cores int) uint64 { return uint64(min(10000, max(1, 100*cores))) }
