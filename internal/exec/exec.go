// Package exec provides the shared subprocess-runner interface used by the
// rest of the codebase to invoke git, gh, and tmux without binding production
// call sites to os/exec directly. Tests inject fakes; production uses Default.
//
// It also owns the constructors for every git and gh subprocess: GitCommand
// (runs git under LC_ALL=C, so the English stderr callers match on is what
// git actually prints) and GhCommand (runs gh with prompts and the update
// notifier disabled). TestNoRawGitGhExec fails on any raw
// exec.Command("git"|"gh", …) in production code outside command.go. tmux
// has its own constructor, session/tmux.Command, enforced the same way.
//
// Kept as a leaf package so every layer — cmd/, session/git/, tools/ — can
// import it without creating a cycle (cmd/ imports session/git/).
package exec

import (
	"os/exec"
	"strings"
)

// Executor runs *exec.Cmd values. Production callers use Default; tests
// substitute a recorder or an in-memory fake.
type Executor interface {
	Run(c *exec.Cmd) error
	Output(c *exec.Cmd) ([]byte, error)
	CombinedOutput(c *exec.Cmd) ([]byte, error)
}

// Default is the production Executor that forwards directly to *exec.Cmd.
type Default struct{}

func (Default) Run(c *exec.Cmd) error                      { return c.Run() }
func (Default) Output(c *exec.Cmd) ([]byte, error)         { return c.Output() }
func (Default) CombinedOutput(c *exec.Cmd) ([]byte, error) { return c.CombinedOutput() }

// ToString renders a command for logs in "argv joined by spaces" form.
func ToString(c *exec.Cmd) string {
	if c == nil {
		return "<nil>"
	}
	return strings.Join(c.Args, " ")
}
