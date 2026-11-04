package sched

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnsupported is what the Windows stubs answer in place of running,
// probing or signalling a job: the runner leans on sessions, process groups
// and flock, which Windows has no counterpart for here.
var ErrUnsupported = errors.New("the work queue isn't available on Windows")

// Runner starts the shim processes that run jobs.
type Runner struct {
	// Shim is the command line before the job dir: {exe, "job-shim"}.
	Shim []string
	// Env is added to the shim process's own environment. It never reaches
	// the job, whose environment comes from its spec.json alone.
	Env []string
}

// NewRunner returns a Runner that starts this process's own executable as
// "job-shim".
func NewRunner() (Runner, error) {
	exe, err := os.Executable()
	if err != nil {
		return Runner{}, fmt.Errorf("finding loom's own executable: %w", err)
	}
	return Runner{Shim: []string{exe, "job-shim"}}, nil
}

// shimEnv is the shim process's environment: this process's own, then r.Env.
// Of two entries for one variable the later wins, as exec.Cmd.Env defines.
func (r Runner) shimEnv() []string {
	return append(os.Environ(), r.Env...)
}
