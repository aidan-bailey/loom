// Command loomdev manages isolated loom dev sandboxes so a dev build can be
// run, driven, and screenshotted from inside loom without touching the host
// loom's tmux sessions or state. See .claude/skills/loom-dev/SKILL.md and
// docs/superpowers/specs/2026-09-16-dev-sandbox-design.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/spf13/cobra"
)

const modulePath = "github.com/aidan-bailey/loom"

type app struct {
	out, errOut io.Writer
	sandbox     string
}

// exitError carries the dev loom's own exit status out of `run`.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("loom exited with status %d", e.code) }

func main() {
	if err := newRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		fmt.Fprintln(os.Stderr, "loomdev:", err)
		os.Exit(1)
	}
}

func newRootCmd(out, errOut io.Writer) *cobra.Command {
	a := &app{out: out, errOut: errOut}
	root := &cobra.Command{
		Use:           "loomdev",
		Short:         "Isolated loom-in-loom dev sandboxes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVarP(&a.sandbox, "sandbox", "s", "",
		"sandbox name (default: leaf of the current git branch)")
	root.AddCommand(a.upCmd(), a.buildCmd(), a.runCmd(), a.startCmd(), a.stopCmd(),
		a.keysCmd(), a.shotCmd(), a.waitCmd(), a.logsCmd(), a.envCmd(), a.lsCmd(), a.downCmd())
	return root
}

// moduleRoot returns the top of the loom checkout loomdev runs in.
func moduleRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("loomdev must run inside a loom checkout: %w", err)
	}
	root := strings.TrimSpace(string(out))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(data), "module "+modulePath+"\n") {
		return "", fmt.Errorf("%s is not a %s checkout", root, modulePath)
	}
	return root, nil
}

func (a *app) open() (*devsandbox.Sandbox, error) {
	name := a.sandbox
	if name == "" {
		out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			return nil, fmt.Errorf("derive the sandbox name from the branch (or pass --sandbox): %w", err)
		}
		name = devsandbox.DefaultName(strings.TrimSpace(string(out)))
	}
	return devsandbox.Open(name)
}

// prepare brings the sandbox up without changing its config, then rebuilds
// it unless skipBuild is set.
func (a *app) prepare(skipBuild bool) (*devsandbox.Sandbox, error) {
	sb, err := a.open()
	if err != nil {
		return nil, err
	}
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	if err := sb.Up(devsandbox.UpOptions{SourceWorktree: root, Warn: a.errOut}); err != nil {
		return nil, err
	}
	if skipBuild {
		if _, err := os.Stat(sb.LoomBin()); err != nil {
			return nil, fmt.Errorf("sandbox %q has no build yet; drop --no-build", sb.Name)
		}
		return sb, nil
	}
	return sb, sb.Build(root)
}
