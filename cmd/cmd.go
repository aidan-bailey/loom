package cmd

import (
	"os/exec"
	"strings"
)

type Executor interface {
	Run(cmd *exec.Cmd) error
	Output(cmd *exec.Cmd) ([]byte, error)
	CombinedOutput(cmd *exec.Cmd) ([]byte, error)
}

type Exec struct{}

func (e Exec) Run(cmd *exec.Cmd) error {
	return cmd.Run()
}

func (e Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	return cmd.Output()
}

func (e Exec) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func MakeExecutor() Executor {
	return Exec{}
}

func ToString(cmd *exec.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	return strings.Join(cmd.Args, " ")
}
