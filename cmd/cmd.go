package cmd

import (
	"os/exec"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// Executor is re-exported from internal/exec so session/git and cmd share
// the same interface without either importing the other (cmd imports
// session/git already).
type Executor = internalexec.Executor

// Exec is the production implementation, aliased from internal/exec.Default.
type Exec = internalexec.Default

// MakeExecutor returns a production Executor.
func MakeExecutor() Executor {
	return internalexec.Default{}
}

// ToString renders a command for logs in "argv joined by spaces" form.
func ToString(cmd *exec.Cmd) string {
	return internalexec.ToString(cmd)
}
