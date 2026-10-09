package cmd_test

import (
	"os/exec"
)

type MockCmdExec struct {
	RunFunc            func(cmd *exec.Cmd) error
	OutputFunc         func(cmd *exec.Cmd) ([]byte, error)
	CombinedOutputFunc func(cmd *exec.Cmd) ([]byte, error)
}

func (e MockCmdExec) Run(cmd *exec.Cmd) error {
	return e.RunFunc(cmd)
}

func (e MockCmdExec) Output(cmd *exec.Cmd) ([]byte, error) {
	return e.OutputFunc(cmd)
}

func (e MockCmdExec) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if e.CombinedOutputFunc != nil {
		return e.CombinedOutputFunc(cmd)
	}
	return e.OutputFunc(cmd)
}

// TmuxSubcommand is the command a tmux argv runs: the first word after the
// global flags tmux.Command puts first (-u, and -L <socket>), or "" when
// there is none. Mocks dispatch on it rather than on argv[1], which is a
// global flag.
func TmuxSubcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		switch argv[i] {
		case "-u":
		case "-L":
			i++ // its socket name
		default:
			return argv[i]
		}
	}
	return ""
}
