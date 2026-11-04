package sched

import (
	"strconv"
	"strings"
)

// jobIDVar names the variable that tells a job its own ID.
const jobIDVar = "LOOM_JOB"

// Hints returns a new environment: env with the parallelism variables it
// lacks added, each set to cpus unless env already sets it (to anything, an
// empty value included), and LOOM_JOB set to id, replacing one a job
// submitted from inside another job inherited. The hints are
// CARGO_BUILD_JOBS=N, RUST_TEST_THREADS=N, MAKEFLAGS=-jN, NIX_BUILD_CORES=N,
// CMAKE_BUILD_PARALLEL_LEVEL=N and GOFLAGS=-p=N. env is left as it is.
func Hints(env []string, cpus int, id JobID) []string {
	n := strconv.Itoa(cpus)
	hints := []struct{ name, value string }{
		{"CARGO_BUILD_JOBS", n},
		{"RUST_TEST_THREADS", n},
		{"MAKEFLAGS", "-j" + n},
		{"NIX_BUILD_CORES", n},
		{"CMAKE_BUILD_PARALLEL_LEVEL", n},
		{"GOFLAGS", "-p=" + n},
	}
	out := make([]string, 0, len(env)+len(hints)+1)
	set := make(map[string]bool, len(env))
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if name == jobIDVar {
			continue
		}
		set[name] = true
		out = append(out, e)
	}
	for _, h := range hints {
		if !set[h.name] {
			out = append(out, h.name+"="+h.value)
		}
	}
	return append(out, jobIDVar+"="+strconv.FormatInt(int64(id), 10))
}
